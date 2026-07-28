import { Create, useForm } from "@refinedev/antd";
import { UserFields } from "./form";

export const UserCreate = () => {
  const { formProps, saveButtonProps } = useForm();
  return (
    <Create saveButtonProps={saveButtonProps}>
      <UserFields formProps={formProps} />
    </Create>
  );
};
