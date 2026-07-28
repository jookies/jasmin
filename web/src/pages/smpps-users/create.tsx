import { Create, useForm } from "@refinedev/antd";
import { SMPPsUserFields } from "./form";

export const SMPPsUserCreate = () => {
  const { formProps, saveButtonProps } = useForm();
  return (
    <Create saveButtonProps={saveButtonProps}>
      <SMPPsUserFields formProps={formProps} />
    </Create>
  );
};
