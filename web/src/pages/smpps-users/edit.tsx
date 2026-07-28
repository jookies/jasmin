import { Edit, useForm } from "@refinedev/antd";
import { SMPPsUserFields } from "./form";

export const SMPPsUserEdit = () => {
  const { formProps, saveButtonProps } = useForm();
  return (
    <Edit saveButtonProps={saveButtonProps}>
      <SMPPsUserFields formProps={formProps} editing />
    </Edit>
  );
};
