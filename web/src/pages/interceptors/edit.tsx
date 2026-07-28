import { Edit, useForm } from "@refinedev/antd";
import { InterceptorFields } from "./form";

export const InterceptorEdit = () => {
  const { formProps, saveButtonProps } = useForm();
  return (
    <Edit saveButtonProps={saveButtonProps}>
      <InterceptorFields formProps={formProps} editing />
    </Edit>
  );
};
